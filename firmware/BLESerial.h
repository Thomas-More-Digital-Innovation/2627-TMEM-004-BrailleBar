#pragma once
#include <Arduino.h>
#include <BLEDevice.h>
#include <BLEServer.h>
#include <BLEUtils.h>
#include <BLE2902.h>

class BLESerialClass : public BLEServerCallbacks, public BLECharacteristicCallbacks {
    typedef std::function<void(uint8_t* data, size_t len)> DataCb;
    typedef std::function<void(bool connected)> StateCb;

   public:
    // запустить
    void begin(const char* name, uint16_t maxTX = 128, uint16_t serviceUUID = 0xFFE0, uint16_t rxUUID = 0xFFE1, uint16_t txUUID = 0xFFE2) {
        _state = false;
        _max_tx = maxTX;

        BLEDevice::init(name);
        BLEDevice::setMTU(_max_tx + 3);

        BLEServer* pServer = BLEDevice::createServer();
        pServer->setCallbacks(this);

        BLEService* pService = pServer->createService(serviceUUID);

        // HM-10 style: apps often read/subscribe on FFE1 too, so it needs notify + CCCD
        _rx = pService->createCharacteristic(rxUUID, BLECharacteristic::PROPERTY_WRITE | BLECharacteristic::PROPERTY_WRITE_NR |
                                                         BLECharacteristic::PROPERTY_READ | BLECharacteristic::PROPERTY_NOTIFY);
        _rx->addDescriptor(new BLE2902());
        _rx->setCallbacks(this);
        _tx = pService->createCharacteristic(txUUID, BLECharacteristic::PROPERTY_NOTIFY);
        _tx->addDescriptor(new BLE2902());
        
        pService->start();

        BLEAdvertising* adv = pServer->getAdvertising();
        adv->addServiceUUID(serviceUUID);
        adv->setScanResponse(false);
        adv->setMinPreferred(0x00);
        adv->start();
    }

    // остановить
    void end() {
        BLEDevice::deinit(true);

        _rx = nullptr;
        _tx = nullptr;
        _state = false;
    }

    // отправить
    void send(const uint8_t* data, size_t len) {
        if (!_tx || !_state || !data) return;

        while (len) {
            size_t tlen = (len < _max_tx) ? len : _max_tx;

            _tx->setValue(data, tlen);
            _tx->notify();
            _rx->setValue(data, tlen);
            _rx->notify();

            yield();

            data += tlen;
            len -= tlen;
        }
    }

    void send(const String& data) {
        send((const uint8_t*)data.c_str(), data.length());
    }

    void send(const char* data) {
        if (!data) return;
        send((const uint8_t*)data, strlen(data));
    }

    // обработчик данных вида f(uint8_t* data, size_t len)
    void onData(DataCb cb) {
        _data_cb = cb;
    }

    // обработчик смены подключения вида f(bool state)
    void onState(StateCb cb) {
        _state_cb = cb;
    }

    // состояние подключения
    bool getState() {
        return _state;
    }

   private:
    BLECharacteristic* _rx = nullptr;
    BLECharacteristic* _tx = nullptr;

    DataCb _data_cb = nullptr;
    StateCb _state_cb = nullptr;

    uint16_t _max_tx = 20;
    bool _state = false;

    void onConnect(BLEServer* ps) override {
        _state = true;
        if (_state_cb) _state_cb(true);
    }

    void onDisconnect(BLEServer* ps) override {
        _state = false;
        if (_state_cb) _state_cb(false);
        ps->getAdvertising()->start();
    }

    void onWrite(BLECharacteristic* ch) override {
        if (ch != _rx) return;
        if (_data_cb) _data_cb(ch->getData(), ch->getLength());
    }
};

extern BLESerialClass BLESerial;